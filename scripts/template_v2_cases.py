"""Synthetic, visibly non-official examples shared by structural and HTTP E2E checks."""
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
PROFILE_DIR=ROOT/'docs/template-profiles/v2'


def cases():
    for path in sorted(PROFILE_DIR.glob('*.json')):
        p=json.loads(path.read_text(encoding='utf-8'))
        values={}
        for f in p['fields']:
            if f['type']=='repeater':
                values[f['key']]=[{i['key']:f"Тест {i['label']} {n}" for i in f['item_fields'] if i.get('required',True)} for n in range(1,4)]
            elif f['type']=='date': values[f['key']]='2026-12-15'
            elif f['type']=='choice': values[f['key']]=f['options'][0]
            elif f['type']=='text_list': values[f['key']]=['Тестовый проект Альфа','Тестовый проект Бета']
            else: values[f['key']]='Тест: '+f['label'].lower()
        values.update(organization='Учебная тестовая организация',signer='Тестов Т. Т.',
                      signer_position='Заместитель Председателя Организации',affected_departments=['Тестовое подразделение по компетенции'])
        # Remove any key not declared in this particular profile.
        values={k:v for k,v in values.items() if k in {f['key'] for f in p['fields']}}
        specific=dict(projects=['«Электронный архив»','«Единый документооборот»'],
                      processes=['учёт документов','хранение документов'],
                      system_full_name='«Учебный архив»',system_short_name='Архив',
                      readiness_document='Протокола приёмочных испытаний',readiness_number='Т-12',readiness_date='2026-12-01',
                      development_document='Техническим заданием',development_date='2026-10-01',development_number='ТЗ-1',
                      commissioning_date='2026-12-15',date_exception='no',change_kind='developed',
                      operating_docs_confirmation='Сведения пользователя: решение от 01.12.2026 № Т-12; файл требует отдельной проверки',
                      group_subject='внедрению электронного архива',purpose='подготовки предложений по внедрению электронного архива',
                      actions='проанализировать процессы и подготовить предложения',result='проект плана мероприятий',
                      addressee='Заместителю Председателя Организации',termination='представления плана мероприятий',
                      leader='начальник отдела ДОУ Тестов А. А.',secretary='специалист отдела ДОУ Тестова Б. Б.',
                      controller='директора департамента ДОУ Тестова В. В.',chair='директор департамента ИТ Тестов Г. Г.',
                      deputy='начальник управления ИТ Тестова Д. Д.',
                      repeal='no',self_control='no',old_act_type='приказ Организации',old_act_date='2026-09-01',old_act_number='Т-9',old_act_name='Об организации работ',
                      start_date='2026-12-01',end_date='2026-12-10',report_deadline='2026-12-15',plan_deadline='2026-12-20',
                      include_source='no',deadline_in_basis='no',independence_required='unknown',personnel_change='yes',access_changed='yes',
                      document_type='Положение',document_name='О ведении учебного архива',effective_date='2027-01-01',transition='',
                      it_department='Департаменту информационных технологий',customer='Департаменту ДОУ',
                      basis='решением руководства Организации от 24.10.2026 № Т-1',
                      module_current_confirmed='no',signer_authority_confirmed='no')
        values.update({k:v for k,v in specific.items() if k in values})
        overrides={
            'working-group':{},
            'project-committee':dict(purpose='координации участников и контроля сроков проектов',basis='решением руководства от 24.10.2026 № Т-1',responsible_unit='Департаментом информационных технологий',old_act_type='распоряжение Организации',old_act_name='О создании проектного комитета'),
            'is-commissioning':dict(purpose='учёту, хранению и поиску электронных документов',responsible='директор Департамента информационных технологий Тестов В. В.'),
            'local-document':dict(purpose='установления порядка ведения учебного архива',responsible_unit='Департаменту ДОУ',location='на внутреннем портале Организации'),
            'records-responsibles':dict(basis='Инструкции по делопроизводству, утверждённой приказом от 24.10.2026 № Т-1',notification_recipient='Департамент ДОУ',coordinator='Департаменту ДОУ'),
            'property-transfer':dict(basis='назначением нового начальника отдела ДОУ',giver='начальнику отдела ДОУ Тестову А. А.',receiver='заместителю начальника отдела ДОУ Тестовой Б. Б.',items='отдела ДОУ: документы по номенклатуре дел и средства доступа',representative='главного специалиста Департамента ДОУ Тестова Г. Г.',transfer_source='Департамента ДОУ',deadline_basis='Положение о передаче дел, пункт 5; срок 15.12.2026',signer_position='Председатель Организации'),
            'internal-audit':dict(basis='годовым планом внутреннего аудита на 2026 год, утверждённым 24.10.2026',unit='Департаменту ДОУ',report_addressee='Председателю Организации',plan_owner='Департаменту ДОУ',access_scope='Доступ предоставляется согласно программе аудита.'),
        }
        values.update(overrides[path.stem])
        yield path.stem,p,values


def source_path(p):
    return ROOT/'data/demo'/p['field_source']['name']
